//! Audited derive of `ShmLayout` for module configuration bodies.
//!
//! This is the only place outside the sys crate that implements the
//! `unsafe` layout trait. The generated impl is sound because the derive
//! accepts only a non-generic `#[repr(C)]` struct with named fields and
//! requires every field type to implement `ShmLayout` itself, which rules
//! out `bool`, enums, references and raw pointers: none of them implement
//! it. Validation and the C-object walk recurse into every field, and the
//! fingerprint mixes every field's offset and fingerprint with the struct's
//! size and alignment.

#![forbid(unsafe_code)]

use proc_macro::TokenStream;
use proc_macro2::TokenStream as TokenStream2;
use quote::{quote, quote_spanned};
use syn::{Data, DeriveInput, Fields, parse_macro_input, spanned::Spanned};

/// Derives `ShmLayout` for a configuration body.
#[proc_macro_derive(ShmLayout)]
pub fn derive_shm_layout(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    match expand(&input) {
        Ok(tokens) => tokens.into(),
        Err(err) => err.to_compile_error().into(),
    }
}

fn is_repr_c(input: &DeriveInput) -> syn::Result<bool> {
    let mut repr_c = false;
    for attr in input.attrs.iter().filter(|a| a.path().is_ident("repr")) {
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("C") {
                repr_c = true;
                Ok(())
            } else if meta.path.is_ident("align") {
                // Alignment changes size and offsets, which the fingerprint
                // covers; the value itself is not needed here.
                let content;
                syn::parenthesized!(content in meta.input);
                let _: syn::LitInt = content.parse()?;
                Ok(())
            } else {
                Err(meta.error("ShmLayout supports only repr(C), optionally with align"))
            }
        })?;
    }
    Ok(repr_c)
}

fn expand(input: &DeriveInput) -> syn::Result<TokenStream2> {
    let name = &input.ident;
    if !input.generics.params.is_empty() {
        return Err(syn::Error::new(
            input.generics.span(),
            "ShmLayout cannot be derived for a generic type",
        ));
    }
    if !is_repr_c(input)? {
        return Err(syn::Error::new(name.span(), "ShmLayout requires #[repr(C)]"));
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
    let shm = quote!(::yanet_sdk::shm);

    let mut fingerprint = Vec::new();
    let mut validate = Vec::new();
    let mut visit = Vec::new();
    let mut bounds = Vec::new();
    for field in &fields.named {
        let ident = field.ident.as_ref().expect("named field");
        let ty = &field.ty;
        bounds.push(quote_spanned! {ty.span()=>
            const _: fn() = || {
                fn field_is_shm_layout<T: #shm::ShmLayout>() {}
                field_is_shm_layout::<#ty>();
            };
        });
        fingerprint.push(quote! {
            hash = #shm::fingerprint_field(
                hash,
                ::core::mem::offset_of!(#name, #ident),
                <#ty as #shm::ShmLayout>::FINGERPRINT,
            );
        });
        validate.push(quote! {
            <#ty as #shm::ShmLayout>::validate(v, addr + ::core::mem::offset_of!(#name, #ident))?;
        });
        visit.push(quote! {
            <#ty as #shm::ShmLayout>::visit_c_objects(addr + ::core::mem::offset_of!(#name, #ident), out);
        });
    }

    Ok(quote! {
        #(#bounds)*

        // SAFETY: a non-generic repr(C) struct whose every field implements
        // the layout trait (checked above) accepts any bit pattern; the
        // generated walks recurse into every field.
        unsafe impl #shm::ShmLayout for #name {
            const FINGERPRINT: u64 = {
                let mut hash = #shm::fingerprint_struct(
                    ::core::mem::size_of::<#name>(),
                    ::core::mem::align_of::<#name>(),
                );
                #(#fingerprint)*
                hash
            };

            fn validate(v: &#shm::Validator<'_>, addr: usize) -> ::core::result::Result<(), #shm::ValidationError> {
                #(#validate)*
                let _ = (v, addr);
                ::core::result::Result::Ok(())
            }

            fn visit_c_objects(addr: usize, out: &mut dyn ::core::ops::FnMut(#shm::CObject)) {
                #(#visit)*
                let _ = (addr, out);
            }
        }
    })
}
