//! `#[derive(ShmLayout)]`.

use proc_macro2::TokenStream;
use quote::{format_ident, quote, quote_spanned};
use syn::{Data, DeriveInput, Fields, GenericParam, Generics, LitInt, spanned::Spanned};

/// Checks the representation: `C`, optionally with alignments, which it
/// returns; the expansion bounds each by the maximum shared-memory alignment.
///
/// The compiler takes the largest of several alignment hints, across one
/// attribute or several, so every one of them is returned.
fn check_repr(input: &DeriveInput) -> syn::Result<Vec<LitInt>> {
    let mut repr_c = false;
    let mut aligns = Vec::new();
    for attr in input.attrs.iter().filter(|a| a.path().is_ident("repr")) {
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("C") {
                repr_c = true;
                Ok(())
            } else if meta.path.is_ident("align") {
                let content;
                syn::parenthesized!(content in meta.input);
                aligns.push(content.parse::<LitInt>()?);
                Ok(())
            } else if meta.path.is_ident("packed") {
                Err(meta.error("ShmLayout rejects repr(packed): fields could be misaligned"))
            } else {
                Err(meta.error("ShmLayout supports only repr(C), optionally with align"))
            }
        })?;
    }
    if repr_c {
        Ok(aligns)
    } else {
        Err(syn::Error::new(input.ident.span(), "ShmLayout requires #[repr(C)]"))
    }
}

/// The struct's generics with every type parameter bound by `Build`, and the
/// parameters as the arguments that name the struct.
///
/// Shared memory holds only `'static` data, so a lifetime is refused.
fn bind_generics(generics: &Generics, p: &TokenStream) -> syn::Result<(Generics, Vec<TokenStream>)> {
    let mut bound = generics.clone();
    let mut args = Vec::new();
    for param in &mut bound.params {
        match param {
            GenericParam::Type(ty) if ty.ident == "__W" => {
                return Err(syn::Error::new(
                    ty.ident.span(),
                    "ShmLayout reserves the parameter name `__W` for the owner of the parts",
                ));
            }
            GenericParam::Type(ty) => {
                ty.bounds.push(syn::parse_quote!(#p::Build));
                let ident = &ty.ident;
                args.push(quote!(#ident));
            }
            GenericParam::Const(konst) if konst.ident == "__W" => {
                return Err(syn::Error::new(
                    konst.ident.span(),
                    "ShmLayout reserves the parameter name `__W` for the owner of the parts",
                ));
            }
            GenericParam::Const(konst) => {
                let ident = &konst.ident;
                args.push(quote!(#ident));
            }
            GenericParam::Lifetime(lifetime) => {
                return Err(syn::Error::new(
                    lifetime.span(),
                    "ShmLayout cannot be derived for a type with a lifetime: shared memory holds only 'static data",
                ));
            }
        }
    }
    Ok((bound, args))
}

pub fn expand(input: &DeriveInput) -> syn::Result<TokenStream> {
    let name = &input.ident;
    let Data::Struct(data) = &input.data else {
        return Err(syn::Error::new(
            name.span(),
            "ShmLayout can only be derived for a struct",
        ));
    };
    let Fields::Named(fields) = &data.fields else {
        return Err(syn::Error::new(name.span(), "ShmLayout requires named fields"));
    };
    if fields.named.is_empty() {
        return Err(syn::Error::new(
            name.span(),
            "ShmLayout requires at least one field; use `()` for an empty configuration",
        ));
    }
    let aligns = check_repr(input)?;
    let sdk = format_ident!("__yanet_sdk_for_{}", name);
    let core = format_ident!("__yanet_core_for_{}", name);
    let p = quote!(#sdk::__private);

    let generic = !input.generics.params.is_empty();
    let (generics, args) = bind_generics(&input.generics, &p)?;
    let (impl_generics, ty_generics, where_clause) = generics.split_for_impl();
    let predicates: Vec<_> = where_clause.iter().flat_map(|w| w.predicates.iter()).collect();
    let ty = quote!(#name #ty_generics);

    let mut bounds = Vec::new();
    let mut parts_fields = Vec::new();
    let mut writes = Vec::new();
    let mut guards = Vec::new();
    let mut frees = Vec::new();
    let mut debug_bounds = Vec::new();
    let mut idents = Vec::new();
    let mut field_idents = Vec::new();
    let mut field_names = Vec::new();
    for (idx, field) in fields.named.iter().enumerate() {
        let ident = field.ident.as_ref().expect("named field");
        let field_ty = &field.ty;
        let vis = &field.vis;
        let binding = format_ident!("__f{}", idx);
        let guard = format_ident!("__g{}", idx);
        bounds.push(quote_spanned! {field_ty.span()=>
            __field_is_build::<#field_ty>();
        });
        parts_fields.push(quote_spanned!(field_ty.span()=> #vis #ident: <#field_ty as #p::Build>::Parts<'b, __W>));
        debug_bounds
            .push(quote_spanned!(field_ty.span()=> <#field_ty as #p::Build>::Parts<'b, __W>: #core::fmt::Debug));
        writes.push(quote_spanned! {field_ty.span()=>
            <#field_ty as #p::Build>::write::<__W>(
                #binding,
                __memory,
                __addr + #core::mem::offset_of!(#ty, #ident),
            )?;
            let #guard = #p::Partial::<#field_ty, __W>::new(
                __memory,
                __addr + #core::mem::offset_of!(#ty, #ident),
                1,
            );
        });
        guards.push(guard);
        frees.push(quote_spanned! {field_ty.span()=>
            <#field_ty as #p::Build>::free::<__W>(
                __addr + #core::mem::offset_of!(#ty, #ident),
                __memory,
            );
        });
        idents.push(quote!(#ident: #binding));
        field_idents.push(ident);
        field_names.push(ident.to_string());
    }
    let drop_msg = format!("ShmLayout: `{name}` needs drop; shared-memory types are never dropped");
    let align_msg = format!("ShmLayout: `{name}` is aligned to more than the block allocator's MAX_ALIGN");
    let vis = &input.vis;
    let parts_name = format_ident!("{}Parts", name);
    let name_str = parts_name.to_string();
    let parts_doc = format!("Parts of [`{name}`]: one per field, each written at the field's place.");

    // The parts take the build's lifetime and owner, then the struct's own
    // parameters.
    let mut parts_generics = generics.clone();
    parts_generics.params.insert(0, syn::parse_quote!(__W: #p::Owner));
    parts_generics.params.insert(0, syn::parse_quote!('b));
    let (parts_impl_generics, parts_ty_generics, _) = parts_generics.split_for_impl();
    let parts_where = &generics.where_clause;

    // A non-generic type is checked once here. A generic one cannot name its
    // parameters in a constant, so every alignment it asks for is checked here,
    // a `Drop` impl clashes with the blanket impl below, and the value's
    // layout is checked again where each instance is written or freed.
    let checks = if generic {
        quote! {
            #(#core::assert!(#aligns <= #p::MAX_ALIGN, #align_msg);)*
            #[allow(dead_code)]
            trait __MustNotImplDrop {}
            #[allow(drop_bounds)]
            impl<__T: #core::ops::Drop> __MustNotImplDrop for __T {}
            impl #impl_generics __MustNotImplDrop for #ty #where_clause {}
        }
    } else {
        quote! {
            #core::assert!(!#core::mem::needs_drop::<#name>(), #drop_msg);
            #core::assert!(#core::mem::align_of::<#name>() <= #p::MAX_ALIGN, #align_msg);
        }
    };
    let instance_checks = generic.then(|| {
        quote! {
            const {
                #core::assert!(!#core::mem::needs_drop::<Self>(), #drop_msg);
                #core::assert!(#core::mem::align_of::<Self>() <= #p::MAX_ALIGN, #align_msg);
            }
        }
    });

    Ok(quote! {
        #[allow(non_snake_case)]
        extern crate yanet_sdk as #sdk;
        #[allow(non_snake_case)]
        extern crate core as #core;

        const _: () = {
            fn __field_is_build<T: #p::Build>() {}
            // Generic over the struct's own parameters, so each field is
            // `Build` for every argument the bounds admit.
            fn __fields #impl_generics () #where_clause {
                #(#bounds)*
            }
            #checks
        };

        // SAFETY: a repr(C) struct, not packed, without drop glue, whose
        // every field is shared-memory data for every argument its bounds
        // admit (checked above), accepts every bit pattern. Each field is
        // aligned to at most 64 by its own layout and every alignment hint
        // of the repr is bounded likewise, so the struct is too. Padding is never
        // accessed as data.
        unsafe impl #impl_generics #p::ShmLayout for #ty #where_clause {}

        #[doc = #parts_doc]
        #vis struct #parts_name #parts_generics #parts_where {
            #(#parts_fields),*
        }

        impl #parts_impl_generics #core::fmt::Debug for #parts_name #parts_ty_generics
        where
            #(#predicates,)*
            #(#debug_bounds,)*
        {
            fn fmt(&self, __f: &mut #core::fmt::Formatter<'_>) -> #core::result::Result<(), #core::fmt::Error> {
                __f.debug_struct(#name_str)
                    #(.field(#field_names, &self.#field_idents))*
                    .finish()
            }
        }

        // SAFETY: every field is written at its own offset from the parts of
        // its own type, each exactly once, and nothing else is written; a
        // refused field returns the fields before it, last first, and the
        // parts after it are dropped; the blocks owned are exactly those of
        // the fields, each freed once.
        unsafe impl #impl_generics #p::Build for #ty #where_clause {
            type Parts<'b, __W: #p::Owner> = #parts_name<'b, __W, #(#args),*>;

            unsafe fn write<__W: #p::Owner>(
                __parts: #parts_name<'_, __W, #(#args),*>,
                __memory: &#p::MemoryContext<'_, __W>,
                __addr: usize,
            ) -> #core::result::Result<(), #p::Refusal> {
                #instance_checks
                let #parts_name { #(#idents),* } = __parts;
                // SAFETY: each field lies in the value at the address, which
                // the caller vouches for, and a guard covers the field just
                // written.
                unsafe {
                    #(#writes)*
                    #(#core::mem::forget(#guards);)*
                }
                #core::result::Result::Ok(())
            }

            unsafe fn free<__W: #p::Owner>(
                __addr: usize,
                __memory: &#p::MemoryContext<'_, __W>,
            ) {
                #instance_checks
                // SAFETY: each field lies in the value at the address, which
                // was written whole (caller contract).
                unsafe {
                    #(#frees)*
                }
            }
        }
    })
}
