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

/// The struct's generics with every type parameter bound by `ShmLayout`.
///
/// Shared memory holds only `'static` data, so a lifetime is refused.
fn bind_generics(generics: &Generics, p: &TokenStream) -> syn::Result<Generics> {
    let mut bound = generics.clone();
    for param in &mut bound.params {
        match param {
            GenericParam::Type(ty) => ty.bounds.push(syn::parse_quote!(#p::ShmLayout)),
            GenericParam::Const(_) => {}
            GenericParam::Lifetime(lifetime) => {
                return Err(syn::Error::new(
                    lifetime.span(),
                    "ShmLayout cannot be derived for a type with a lifetime: shared memory holds only 'static data",
                ));
            }
        }
    }
    Ok(bound)
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
    let generics = bind_generics(&input.generics, &p)?;
    let (impl_generics, ty_generics, where_clause) = generics.split_for_impl();
    let ty = quote!(#name #ty_generics);

    let mut bounds = Vec::new();
    let mut layout_fields = Vec::new();
    for field in &fields.named {
        let ident = field.ident.as_ref().expect("named field");
        let field_ty = &field.ty;
        bounds.push(quote_spanned! {field_ty.span()=>
            __field_is_shm_layout::<#field_ty>();
        });
        let layout_name = ident.to_string();
        layout_fields.push(quote_spanned! {field_ty.span()=>
            #p::LayoutField {
                name: #layout_name,
                offset: #core::mem::offset_of!(#ty, #ident),
                ty: <#field_ty as #p::ShmLayout>::LAYOUT,
            }
        });
    }
    let drop_msg = format!("ShmLayout: `{name}` needs drop; shared-memory types are never dropped");
    let align_msg = format!("ShmLayout: `{name}` is aligned to more than the block allocator's MAX_ALIGN");

    // A non-generic type is checked once here. A generic one cannot name its
    // parameters in a constant, so every alignment it asks for is checked here
    // and a `Drop` impl clashes with the blanket impl below.
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

    Ok(quote! {
        #[allow(non_snake_case)]
        extern crate yanet_sdk as #sdk;
        #[allow(non_snake_case)]
        extern crate core as #core;

        const _: () = {
            fn __field_is_shm_layout<T: #p::ShmLayout>() {}
            // Generic over the struct's own parameters, so each field is
            // `ShmLayout` for every argument the bounds admit.
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
        unsafe impl #impl_generics #p::ShmLayout for #ty #where_clause {
            const LAYOUT: &'static #p::LayoutNode = &#p::LayoutNode::of::<Self>(#p::LayoutKind::Struct {
                name: #core::concat!(#core::module_path!(), "::", #core::stringify!(#name)),
                fields: &[#(#layout_fields),*],
            });
        }
    })
}
