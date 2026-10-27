//! `#[derive(ShmLayout)]`.

use proc_macro2::TokenStream;
use quote::{format_ident, quote, quote_spanned};
use syn::{Data, DeriveInput, Fields, spanned::Spanned};

/// Checks the representation: `C`, optionally with an alignment; the
/// expansion bounds the alignment by `MAX_ALIGN`.
fn check_repr(input: &DeriveInput) -> syn::Result<()> {
    let mut repr_c = false;
    for attr in input.attrs.iter().filter(|a| a.path().is_ident("repr")) {
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("C") {
                repr_c = true;
                Ok(())
            } else if meta.path.is_ident("align") {
                let content;
                syn::parenthesized!(content in meta.input);
                content.parse::<syn::LitInt>()?;
                Ok(())
            } else if meta.path.is_ident("packed") {
                Err(meta.error("ShmLayout rejects repr(packed): fields could be misaligned"))
            } else {
                Err(meta.error("ShmLayout supports only repr(C), optionally with align"))
            }
        })?;
    }
    if repr_c {
        Ok(())
    } else {
        Err(syn::Error::new(input.ident.span(), "ShmLayout requires #[repr(C)]"))
    }
}

pub fn expand(input: &DeriveInput) -> syn::Result<TokenStream> {
    let name = &input.ident;
    if !input.generics.params.is_empty() || input.generics.where_clause.is_some() {
        return Err(syn::Error::new(
            input.generics.span(),
            "ShmLayout cannot be derived for a generic type",
        ));
    }
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
    check_repr(input)?;
    let p = quote!(::yanet_sdk::__private);

    let mut bounds = Vec::new();
    let mut sizes = Vec::new();
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
        let ty = &field.ty;
        let vis = &field.vis;
        let binding = format_ident!("__f{}", idx);
        let guard = format_ident!("__g{}", idx);
        bounds.push(quote_spanned! {ty.span()=>
            __field_is_build::<#ty>();
        });
        sizes.push(quote!(+ ::core::mem::size_of::<#ty>()));
        parts_fields.push(quote_spanned!(ty.span()=> #vis #ident: <#ty as #p::Build>::Parts<'b, __W>));
        debug_bounds.push(quote_spanned!(ty.span()=> <#ty as #p::Build>::Parts<'b, __W>: ::core::fmt::Debug));
        writes.push(quote_spanned! {ty.span()=>
            <#ty as #p::Build>::write::<__W>(
                #binding,
                __memory,
                __addr + ::core::mem::offset_of!(#name, #ident),
            )?;
            let #guard = #p::Partial::<#ty, __W>::new(
                __memory,
                __addr + ::core::mem::offset_of!(#name, #ident),
                1,
            );
        });
        guards.push(guard);
        frees.push(quote_spanned! {ty.span()=>
            <#ty as #p::Build>::free::<__W>(
                __addr + ::core::mem::offset_of!(#name, #ident),
                __memory,
            );
        });
        idents.push(quote!(#ident: #binding));
        field_idents.push(ident);
        field_names.push(ident.to_string());
    }
    let padding_msg = format!("ShmLayout: `{name}` has implicit padding; add explicit padding fields");
    let drop_msg = format!("ShmLayout: `{name}` needs drop; shared-memory types are never dropped");
    let align_msg = format!("ShmLayout: `{name}` is aligned to more than the block allocator's MAX_ALIGN");
    let vis = &input.vis;
    let parts_name = format_ident!("{}Parts", name);
    let name_str = parts_name.to_string();
    let parts_doc = format!("Parts of [`{name}`]: one per field, each written at the field's place.");

    Ok(quote! {
        const _: () = {
            fn __field_is_build<T: #p::Build>() {}
            fn __fields() {
                #(#bounds)*
            }
            ::core::assert!(
                ::core::mem::size_of::<#name>() == 0 #(#sizes)*,
                #padding_msg,
            );
            ::core::assert!(!::core::mem::needs_drop::<#name>(), #drop_msg);
            ::core::assert!(::core::mem::align_of::<#name>() <= #p::MAX_ALIGN, #align_msg);
        };

        // SAFETY: a non-generic repr(C) struct, not packed, aligned to at
        // most 64, without padding (size equals the sum of its fields) or
        // drop glue, whose every field is ShmLayout (checked above), accepts
        // every bit pattern.
        unsafe impl #p::ShmLayout for #name {}

        #[doc = #parts_doc]
        #vis struct #parts_name<'b, __W: #p::Owner> {
            #(#parts_fields),*
        }

        impl<'b, __W: #p::Owner> ::core::fmt::Debug for #parts_name<'b, __W>
        where
            #(#debug_bounds,)*
        {
            fn fmt(&self, __f: &mut ::core::fmt::Formatter<'_>) -> ::core::result::Result<(), ::core::fmt::Error> {
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
        unsafe impl #p::Build for #name {
            type Parts<'b, __W: #p::Owner> = #parts_name<'b, __W>;

            unsafe fn write<__W: #p::Owner>(
                __parts: #parts_name<'_, __W>,
                __memory: &#p::MemoryContext<'_, __W>,
                __addr: usize,
            ) -> ::core::result::Result<(), #p::Refusal> {
                let #parts_name { #(#idents),* } = __parts;
                // SAFETY: each field lies in the value at the address, which
                // the caller vouches for, and a guard covers the field just
                // written.
                unsafe {
                    #(#writes)*
                    #(::core::mem::forget(#guards);)*
                }
                ::core::result::Result::Ok(())
            }

            unsafe fn free<__W: #p::Owner>(
                __addr: usize,
                __memory: &#p::MemoryContext<'_, __W>,
            ) {
                // SAFETY: each field lies in the value at the address, which
                // was written whole (caller contract).
                unsafe {
                    #(#frees)*
                }
            }
        }
    })
}
